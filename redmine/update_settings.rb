require 'yaml'
require 'optparse'

options = {}
OptionParser.new do |opts|
  opts.banner = "Usage: update_settings.rb [options]"
  opts.on("--hostname HOSTNAME", "Set the hostname") do |value|
    options[:hostname] = value
  end
end.parse!

file_path = 'config/settings.yml'
settings = YAML.load_file(file_path)

# General
## Host name and path
settings['host_name']['default'] = options[:hostname] + ':9441'
## Protocol
settings['protocol']['default'] = 'https'
## Text formatting
settings['text_formatting']['default'] = 'common_mark'

# Display
## Force default language for anonymous users
settings['force_default_language_for_anonymous']['default'] = 1
## Force default language for logged-in users
settings['force_default_language_for_loggedin']['default'] = 1

# Authentication
## Authentication required
settings['login_required']['default'] = 1
## Self-registration
settings['self_registration']['default'] = '0'
## Allow password reset via email
settings['lost_password']['default'] = 0
## Two-factor authentication
settings['twofa']['default'] = 0

# Projects
## New projects are public by default
settings['default_projects_public']['default'] = 0

# Users
## Maximum number of additional email addresses
settings['max_additional_emails']['default'] = 0
## Allowed email domains
settings['email_domains_allowed']['default'] = options[:hostname]
## Allow users to delete their own account
settings['unsubscribe']['default'] = 0

# Issue tracking
## Allow cross-project issue relations
settings['cross_project_issue_relations']['default'] = 1
## Link issues on copy
settings['link_copied_issue']['default'] = 'no'
## Allow cross-project subtasks
settings['cross_project_subtasks']['default'] = 'system'
## Use current date as start date for new issues
settings['default_issue_start_date_to_creation_date']['default'] = 0
## Display subprojects issues on main projects by default
settings['display_subprojects_issues']['default'] = 0

# Email notifications
## Emission email address
settings['mail_from']['default'] = 'redmine@' + options[:hostname]

File.open(file_path, 'w') { |file| file.write(settings.to_yaml) }
puts "Updated #{file_path} successfully."
